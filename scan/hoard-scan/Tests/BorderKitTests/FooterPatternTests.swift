import Foundation
import Testing

@testable import BorderKit

private func firstGroup(_ re: NSRegularExpression, _ s: String) -> String? {
    let range = NSRange(s.startIndex..., in: s)
    guard let m = re.firstMatch(in: s, range: range),
          m.numberOfRanges > 1,
          let r = Range(m.range(at: 1), in: s) else { return nil }
    return String(s[r])
}

@Test("the copyright range captures the year that dates the printing")
func copyrightRangeCaptures() {
    #expect(firstGroup(copyrightYearRE, "© 1993-2003 Wizards of the Coast, Inc.") == "2003")
    #expect(firstGroup(copyrightYearRE, "© 1993 2003 Wizards") == "2003")
}

@Test("the collector pair captures number over total")
func collectorPairCaptures() {
    #expect(firstGroup(collectorPairRE, "95/350") == "95")
    #expect(firstGroup(collectorPairRE, "no numbers here") == nil)
}

@Test("a lone year still reads on the frames that print no range")
func loneYearCaptures() {
    #expect(firstGroup(copyrightLoneYearRE, "© 1995 Wizards") == "1995")
}

@Test("the never-match fallback truly never matches")
func fallbackNeverMatches() {
    let re = footerPattern("(this[is(not(a(pattern")
    let s = "© 1993-2003 Wizards 95/350"
    #expect(re.firstMatch(in: s, range: NSRange(s.startIndex..., in: s)) == nil)
}
