import Foundation

public func editDistance(_ a: String, _ b: String) -> Int {
    let x = Array(a), y = Array(b)
    if x.isEmpty { return y.count }
    if y.isEmpty { return x.count }
    var prev = Array(0...y.count)
    var cur = [Int](repeating: 0, count: y.count + 1)
    for i in 1...x.count {
        cur[0] = i
        for j in 1...y.count {
            let sub = prev[j - 1] + (x[i - 1] == y[j - 1] ? 0 : 1)
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, sub)
        }
        swap(&prev, &cur)
    }
    return prev[y.count]
}

public func copyrightFurniture(_ s: String) -> Bool {
    let tokens = s.lowercased().split(whereSeparator: { !$0.isLetter && !$0.isNumber })
    let brand = tokens.contains("coast") || tokens.contains(where: { $0.hasPrefix("wizar") })
    guard brand else { return false }
    if s.contains("©") || s.contains("™") { return true }
    if tokens.contains(where: { ["inc", "ine", "in", "ir", "lnc"].contains($0) }) { return true }
    let ascii = asciify(s)
    if group(copyrightYearRE, ascii) != nil { return true }
    if group(collectorPairRE, ascii) != nil { return true }
    if let y = group(copyrightLoneYearRE, ascii), let n = Int(y), n >= 1993, n <= 2035 {
        return true
    }
    return false
}

public func artistCredit(_ s: String) -> Bool {
    let words = s.split(whereSeparator: { $0.isWhitespace }).map(String.init)
    guard words.count == 3 else { return false }
    guard let last = words[0].last, last == "." || last == "," else { return false }
    let head = words[0].lowercased().filter { $0.isLetter }
    guard head.count >= 3, head.count <= 6 else { return false }
    guard editDistance(head, "illus") <= 4 else { return false }
    return words.dropFirst().allSatisfy { $0.first?.isUppercase == true }
}
