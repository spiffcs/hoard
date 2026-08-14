import Foundation

public let copyrightYearRE = footerPattern(
    #"(?:19|20)\d{2}\s*[-–—]?\s*((?:19|20)\d{2})"#)
public let collectorPairRE = footerPattern(
    #"(\d{1,5})\s*/\s*(\d{1,5})"#)
public let copyrightLoneYearRE = footerPattern(
    #"\b((?:19|20)\d{2})\b"#)

func footerPattern(_ pattern: String) -> NSRegularExpression {
    (try? NSRegularExpression(pattern: pattern))
        ?? (try? NSRegularExpression(pattern: "(?!)"))
        ?? NSRegularExpression()
}
