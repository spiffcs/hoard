import Foundation

public struct Printing: Equatable, Sendable {
    public var number = ""
    public var total: Int?
    public var setCode = ""
    public var rarity = ""
    public var language = ""
    public var finish = ""
    public var finishSource = ""
    public var year: Int?
    public var yearFrom: Int?
    public var numberSource: NumberSource = .none

    public enum NumberSource: String, Equatable, Sendable {
        case none, ownRow, copyrightRow
    }

    public var isEmpty: Bool {
        number.isEmpty && setCode.isEmpty && year == nil
    }
}

public func readPrinting(bandLines lines: [String]) -> Printing {
    var out = Printing()
    var ownRowIsBare = false

    for line in lines {
        if looksLikeCompanyRow(line) || line.contains("©") {
            var consumed = false
            if let (from, to) = years(in: line) {
                consumed = true
                if out.year == nil || to > out.year! {
                    out.year = to
                    out.yearFrom = from
                }
            }
            let ownRowHolds = out.numberSource == .ownRow && !ownRowIsBare
            if !ownRowHolds, let pair = collectorPair(in: line) {
                out.number = pair.number
                out.total = pair.total
                out.numberSource = .copyrightRow
                ownRowIsBare = false
                consumed = true
            } else if out.numberSource == .none || ownRowIsBare,
                      let n = trailingNumber(in: line) {
                out.number = n
                out.numberSource = .copyrightRow
                ownRowIsBare = false
                consumed = true
            }
            if consumed { continue }
        }

        if let set = setRow(in: line) {
            out.setCode = set.code
            out.language = set.language
            if out.finish.isEmpty, !set.finish.isEmpty {
                out.finish = set.finish
                out.finishSource = "separator"
            }
            continue
        }

        if let n = ownNumberRow(line) {
            if n.bare && out.numberSource == .copyrightRow {
                if out.rarity.isEmpty { out.rarity = n.rarity }
                continue
            }
            out.number = n.number
            out.rarity = n.rarity
            if let total = n.total { out.total = total }
            out.numberSource = .ownRow
            ownRowIsBare = n.bare
        }
    }
    return out
}

func years(in line: String) -> (from: Int?, to: Int)? {
    guard looksLikeCompanyRow(line) || line.contains("©") else { return nil }
    let tokens = line.split { !$0.isLetter && !$0.isNumber }
    let found = tokens.compactMap { year(from: $0) }
    guard let last = found.last else { return nil }
    return (found.count > 1 ? found.first : nil, last)
}

func collectorPair(in line: String) -> (number: String, total: Int)? {
    let parts = line.split(separator: "/")
    guard parts.count == 2 else { return nil }
    guard let numTok = parts[0].split(separator: " ").last,
          let number = digitsOnly(numTok), number.count <= 4,
          let totalTok = parts[1].split(separator: " ").first,
          let totalStr = digitsOnly(totalTok), let total = Int(totalStr),
          total > 0, total < 10000
    else { return nil }
    return (stripLeadingZeros(number), total)
}

func finishFromSeparator(_ sep: String) -> String {
    if sep.isEmpty { return "" }
    if sep.contains(where: { "★✦✧✶*+".contains($0) }) { return "foil" }
    if sep.contains(where: { "•·∙.,:;|/\\―—–-".contains($0) }) { return "nonfoil" }
    return ""
}

func alphanumericTokens(_ line: String) -> [(text: String, start: String.Index, end: String.Index)] {
    var out: [(String, String.Index, String.Index)] = []
    var i = line.startIndex
    while i < line.endIndex {
        guard line[i].isLetter || line[i].isNumber else {
            i = line.index(after: i)
            continue
        }
        let start = i
        while i < line.endIndex, line[i].isLetter || line[i].isNumber {
            i = line.index(after: i)
        }
        out.append((String(line[start..<i]), start, i))
    }
    return out
}

func trailingNumber(in line: String) -> String? {
    let words = line.split(separator: " ")
    guard let last = words.last, let digits = digitsOnly(last) else { return nil }
    guard (1...4).contains(digits.count) else { return nil }
    if let n = Int(digits), plausibleYears.contains(n) { return nil }
    guard words.count >= 2 else { return nil }
    let prev = String(words[words.count - 2]).lowercased().filter { $0.isLetter }
    guard editDistance(prev, "coast") <= 1 || editDistance(prev, "inc") <= 1
            || editDistance(prev, "wizards") <= 2
    else { return nil }
    let stripped = String(digits.drop(while: { $0 == "0" }))
    return stripped.isEmpty ? nil : stripped
}

func setRow(in line: String) -> (code: String, language: String, finish: String)? {
    let tokens = alphanumericTokens(line)
    guard tokens.count >= 2 else { return nil }
    let code = tokens[0].text.uppercased()
    guard (3...5).contains(code.count), code.allSatisfy({ $0.isLetter || $0.isNumber }),
          code.contains(where: { $0.isLetter })
    else { return nil }
    for (offset, tok) in tokens.dropFirst().prefix(2).enumerated() {
        guard tok.text.filter({ $0.isLetter }).allSatisfy({ $0.isUppercase })
        else { continue }
        if offset == 1, tokens[1].text.count > 2 { return nil }
        let lang = tok.text.uppercased().filter { $0.isLetter }
        if lang.count == 2, knownLanguages.contains(lang) {
            let sep = String(line[tokens[0].end..<tok.start])
                .trimmingCharacters(in: .whitespaces)
            return (code, lang, finishFromSeparator(sep))
        }
        if (3...4).contains(lang.count) {
            let head = String(lang.prefix(2))
            if knownLanguages.contains(head) {
                let sep = String(line[tokens[0].end..<tok.start])
                    .trimmingCharacters(in: .whitespaces)
                return (code, head, finishFromSeparator(sep))
            }
        }
        if lang.count == 3, let first = lang.first, "KXAT".contains(first),
           knownLanguages.contains(String(lang.dropFirst())) {
            return (code, String(lang.dropFirst()), "foil")
        }
    }
    return nil
}

let knownLanguages: Set<String> = [
    "EN", "DE", "FR", "IT", "ES", "PT", "JA", "KO", "RU", "ZH", "CS", "CT", "PH",
]

func ownNumberRow(_ line: String) -> (number: String, rarity: String, total: Int?, bare: Bool)? {
    let tokens = line.split(separator: " ").map(String.init)
        .filter { !$0.isEmpty }
    guard let first = tokens.first else { return nil }

    if first.contains("/") {
        let halves = first.split(separator: "/")
        guard halves.count == 2,
              let n = digitsOnly(halves[0]), n.count <= 4,
              let t = digitsOnly(halves[1]), let total = Int(t), total > 0
        else { return nil }
        let trailing = tokens.count > 1
            ? tokens[1].uppercased().filter { $0.isLetter } : ""
        let hasRarity = trailing.count == 1 && knownRarities.contains(trailing)
        guard hasRarity || total >= 100 else { return nil }
        return (stripLeadingZeros(n), hasRarity ? trailing : "", total, false)
    }

    switch tokens.count {
    case 1:
        guard !tokens[0].contains(where: { currencyGlyphs.contains($0) }),
              let d = digitsOnly(tokens[0]), (2...4).contains(d.count)
        else { return nil }
        return (stripLeadingZeros(d), "", nil, true)
    case 2:
        let lead = tokens[0].uppercased().filter { $0.isLetter }
        guard lead.count == 1, knownRarities.contains(lead),
              let d = digitsOnly(tokens[1]), (2...4).contains(d.count)
        else { return nil }
        return (stripLeadingZeros(d), lead, nil, d.count < 3)
    default:
        return nil
    }
}

let currencyGlyphs: Set<Character> = ["$", "£", "€", "¥", "₹"]

let knownRarities: Set<String> = ["C", "U", "R", "M", "S", "T", "L", "P"]

func stripLeadingZeros(_ s: String) -> String {
    let trimmed = String(s.drop { $0 == "0" })
    return trimmed.isEmpty ? "0" : trimmed
}
