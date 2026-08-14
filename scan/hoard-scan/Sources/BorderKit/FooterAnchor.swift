import Foundation

func illusToken(_ s: String) -> Bool {
    guard let first = s.split(whereSeparator: { $0.isWhitespace }).first else { return false }
    guard let last = first.last, last == "." || last == ":" || last == "," else { return false }
    let head = first.lowercased().filter { $0.isLetter }
    guard head.count >= 3, head.count <= 6 else { return false }
    return editDistance(head, "illus") <= 2
}

enum AnchorKind: String {
    case copyright
    case credit
}

func personalNameLine(_ s: String) -> Bool {
    let words = s.split(whereSeparator: { $0.isWhitespace }).map(String.init)
    guard words.count == 2 || words.count == 3 else { return false }
    return words.allSatisfy { w in
        guard let f = w.first, f.isUppercase else { return false }
        return w.dropFirst().allSatisfy { $0.isLetter || $0 == "." || $0 == "'" || $0 == "-" }
    }
}

func footerAnchor(_ lines: [Line]) -> (line: Line, kind: AnchorKind)? {
    let proven = lines.compactMap { line -> (Line, AnchorKind)? in
        if copyrightFurniture(line.text) { return (line, .copyright) }
        if artistCredit(line.text) || illusToken(line.text) { return (line, .credit) }
        return nil
    }
    if let best = proven.min(by: { $0.0.box.midY < $1.0.box.midY }) { return best }

    return positionalCredit(lines).map { ($0, .credit) }
}

func positionalCredit(_ lines: [Line]) -> Line? {
    guard lines.count >= 3 else { return nil }
    let ys = lines.map { $0.box.midY }
    guard let low = ys.min(), let high = ys.max(), high - low > 0.05 else { return nil }
    let foot = low + (high - low) * 0.25
    let candidates = lines.filter { $0.box.midY <= foot && personalNameLine($0.text) }
    guard let credit = candidates.min(by: { $0.box.midY < $1.box.midY }) else { return nil }
    let above = lines.filter { $0.box.midY > credit.box.midY + 0.02 }
    guard above.count >= 2 else { return nil }
    return credit
}
