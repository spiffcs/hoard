import Foundation

private let digitLookalikes: [Character: Character] = [
    "O": "0", "o": "0", "D": "0", "Q": "0",
    "l": "1", "I": "1", "i": "1", "|": "1",
    "S": "5", "s": "5",
    "B": "8",
    "Z": "2", "z": "2",
    "G": "6",
]

func digitsOnly(_ s: some StringProtocol) -> String? {
    var out = ""
    var real = 0
    var dropped = 0
    var considered = 0
    for ch in s {
        if ch == " " || ch == "," || ch == "." { continue }
        considered += 1
        if ch.isNumber {
            out.append(ch)
            real += 1
        } else if let folded = digitLookalikes[ch] {
            out.append(folded)
        } else {
            dropped += 1
        }
    }
    guard !out.isEmpty, dropped <= 1, real * 2 >= considered else { return nil }
    return out
}

func squashed(_ s: some StringProtocol) -> String {
    String(s.lowercased().filter { $0.isLetter || $0.isNumber })
}

func looksLikeCompanyRow(_ line: String) -> Bool {
    let s = squashed(line)
    if s.contains("coast") || s.contains("wizard") || s.contains("wizard5") {
        return true
    }
    return line.split(whereSeparator: { !$0.isLetter }).contains { tok in
        let t = tok.lowercased()
        return (6...8).contains(t.count) && editDistance(t, "wizards") <= 2
    }
}

func editDistance(_ a: some StringProtocol, _ b: some StringProtocol) -> Int {
    let x = Array(a), y = Array(b)
    if x.isEmpty { return y.count }
    if y.isEmpty { return x.count }
    var prev = Array(0...y.count)
    var cur = [Int](repeating: 0, count: y.count + 1)
    for i in 1...x.count {
        cur[0] = i
        for j in 1...y.count {
            cur[j] = x[i - 1] == y[j - 1]
                ? prev[j - 1]
                : 1 + min(prev[j - 1], prev[j], cur[j - 1])
        }
        swap(&prev, &cur)
    }
    return prev[y.count]
}

func looksLikeIllustratorRow(_ line: String) -> Bool {
    let s = squashed(line)
    return s.hasPrefix("illus") || s.hasPrefix("ilus") || s.hasPrefix("fllus")
        || s.hasPrefix("flus") || s.hasPrefix("lllus")
}

let plausibleYears = 1993...2035

func year(from token: some StringProtocol) -> Int? {
    guard let digits = digitsOnly(token), digits.count == 4,
          let n = Int(digits), plausibleYears.contains(n)
    else { return nil }
    return n
}
