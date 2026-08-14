import CoreGraphics

let cardAspect: CGFloat = 63.0 / 88.0

public enum CardLayout {
    static let footerV: CGFloat = 0.9375
    static let creditV: CGFloat = 0.9212
    static let titleV: CGFloat = 0.0625
    static let borderV: CGFloat = 0.039
    static let ringDepth: CGFloat = 0.45
    static let footerGlyphV: CGFloat = 0.0174
    static let innerV: CGFloat = 0.950
    static let copyrightLeftU: CGFloat = 0.086
    static let creditLeftU: CGFloat = 0.097

    static func leftU(kind: AnchorKind, prefix: LinePrefix,
                      frame: FrameEvidence) -> CGFloat? {
        let year = frame.year
        if year == 0 || year < 1998 {
            switch prefix {
            case .copyrightGlyph, .trademark: return 0.086
            case .illus: return 0.097
            case .year: return 0.102
            }
        }
        if year <= 2002 {
            switch prefix {
            case .trademark: return 0.233
            case .year: return 0.260
            case .copyrightGlyph, .illus: return nil
            }
        }
        if frame.isM15 {
            switch prefix {
            case .trademark: return kind == .copyright ? 0.593 : nil
            case .copyrightGlyph: return kind == .copyright ? 0.596 : nil
            case .year, .illus: return nil
            }
        }
        switch prefix {
        case .trademark: return kind == .copyright ? 0.079 : nil
        case .copyrightGlyph, .year, .illus: return nil
        }
    }
    static let symbolU: CGFloat = 0.872
    static let symbolV: CGFloat = 0.584

    public static let sparkleU: CGFloat = 0.205
    public static let sparkleV: CGFloat = 0.889
}

public struct AnchorMeasurement: Sendable {
    public let kind: String
    public let prefix: String
    public let leftU: CGFloat
    public let text: String
}

public func measureAnchor(_ lines: [Line], year: Int) -> AnchorMeasurement? {
    guard let anchor = footerAnchor(lines) else { return nil }
    guard let prefix = lineOpener(anchor.line.text, kind: anchor.kind) else { return nil }
    let name: String
    switch prefix {
    case .trademark: name = "trademark"
    case .copyrightGlyph: name = "copyrightGlyph"
    case .year: name = "year"
    case .illus: name = "illus"
    }
    return AnchorMeasurement(kind: anchor.kind.rawValue, prefix: name,
                             leftU: anchor.line.box.minX, text: anchor.line.text)
}

public struct FrameEvidence: Sendable {
    public let year: Int
    public let hasSetCode: Bool
    public let numberOnOwnRow: Bool

    public init(year: Int, hasSetCode: Bool = false, numberOnOwnRow: Bool = false) {
        self.year = year
        self.hasSetCode = hasSetCode
        self.numberOnOwnRow = numberOnOwnRow
    }

    public var isM15: Bool {
        if year >= 2015 { return true }
        if year == 2014 { return hasSetCode || numberOnOwnRow }
        return false
    }
}
