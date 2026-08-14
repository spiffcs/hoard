import BorderKit
import CardKit
import CoreGraphics
import Foundation
import ImageIO

enum CorpusCrop {
    static let u0: CGFloat = 0
    static let u1: CGFloat = 284.0 / 630.0
    static let v0: CGFloat = 634.0 / 880.0
    static let v1: CGFloat = 1.0
}

struct CorpusCard {
    let width: Int, height: Int
    private let data: [UInt8]

    init?(_ url: URL) {
        guard let src = CGImageSourceCreateWithURL(url as CFURL, nil),
              let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) else { return nil }
        let w = cg.width, h = cg.height
        guard w > 0, h > 0 else { return nil }
        var buf = [UInt8](repeating: 0, count: w * h * 4)
        let ok = buf.withUnsafeMutableBytes { raw -> Bool in
            guard let ctx = CGContext(
                data: raw.baseAddress, width: w, height: h, bitsPerComponent: 8,
                bytesPerRow: w * 4, space: CGColorSpaceCreateDeviceRGB(),
                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
            return true
        }
        guard ok else { return nil }
        width = w; height = h; data = buf
    }

    private func rgb(_ u: CGFloat, _ v: CGFloat) -> (CGFloat, CGFloat, CGFloat)? {
        let x = Int((u - CorpusCrop.u0) / (CorpusCrop.u1 - CorpusCrop.u0) * CGFloat(width))
        let y = Int((v - CorpusCrop.v0) / (CorpusCrop.v1 - CorpusCrop.v0) * CGFloat(height))
        guard x >= 0, x < width, y >= 0, y < height else { return nil }
        let o = (y * width + x) * 4
        return (CGFloat(data[o]) / 255, CGFloat(data[o + 1]) / 255, CGFloat(data[o + 2]) / 255)
    }

    var sampler: CardSampler {
        { u, v in
            guard let c = rgb(u, v) else { return nil }
            return 0.2126 * c.0 + 0.7152 * c.1 + 0.0722 * c.2
        }
    }

    var chromaSampler: CardSampler {
        { u, v in
            guard let c = rgb(u, v) else { return nil }
            return c.2 - c.0
        }
    }
}

struct CorpusRow {
    let id, session, frame, era, finish, physical, name: String
}

func readLabels(_ path: String) -> [CorpusRow] {
    guard let text = try? String(contentsOfFile: path, encoding: .utf8) else {
        die("no labels at \(path)")
    }
    return text.split(whereSeparator: \.isNewline).dropFirst().compactMap { line in
        let f = line.split(separator: "\t", omittingEmptySubsequences: false)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
        guard f.count >= 7 else { return nil }
        return CorpusRow(id: f[0], session: f[1], frame: f[2], era: f[3],
                         finish: f[4], physical: f[5], name: f[6])
    }
}

func alignedPatch(_ card: CorpusCard, _ template: [CGFloat]) -> [CGFloat]? {
    let sample = card.sampler
    let cellU = SparkleGate.searchU / CGFloat(SparkleGate.searchCellsU)
    let cellV = SparkleGate.searchV / CGFloat(SparkleGate.searchCellsV)
    var best = -CGFloat.infinity
    var bestI = 0, bestJ = 0
    for i in -SparkleGate.searchCellsU...SparkleGate.searchCellsU {
        for j in -SparkleGate.searchCellsV...SparkleGate.searchCellsV {
            guard let p = sparklePatch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV,
                                       step: 1, sample),
                  let n = sparkleNormalise(p) else { continue }
            let s = sparkleCorrelate(n, template)
            if s > best { best = s; bestI = i; bestJ = j }
        }
    }
    guard best > -CGFloat.infinity else { return nil }
    return sparklePatch(du: CGFloat(bestI) * cellU, dv: CGFloat(bestJ) * cellV, step: 1, sample)
}

func sparkleFit(dir: String, out: String, onlySession: String?, chroma: Bool = false) -> Never {
    let labels = readLabels("\(dir)/labels.tsv")
    let foils = labels.filter {
        $0.frame == "retro" && $0.finish == "foil"
            && (onlySession == nil || $0.session == onlySession!)
    }
    guard !foils.isEmpty else { die("no foil rows in \(dir)/labels.tsv") }
    let cards = foils.compactMap { row -> CorpusCard? in
        CorpusCard(URL(fileURLWithPath: "\(dir)/cards/\(row.id).png"))
    }
    guard cards.count == foils.count else {
        die("read \(cards.count) of \(foils.count) foil crops from \(dir)/cards")
    }

    let cells = SparkleTemplate.cols * SparkleTemplate.rows
    var template = [CGFloat](repeating: 0, count: cells)

    var acc = [CGFloat](repeating: 0, count: cells)
    var used = 0
    for card in cards {
        let sample = chroma ? card.chromaSampler : card.sampler
        guard let p = sparklePatch(du: 0, dv: 0, step: 1, sample),
              let n = sparkleNormalise(p) else { continue }
        for i in 0..<cells { acc[i] += n[i] }
        used += 1
    }
    guard used > 0, let seeded = sparkleNormalise(acc.map { $0 / CGFloat(used) }) else {
        die("could not seed a template")
    }
    template = seeded

    for pass in 1...3 {
        acc = [CGFloat](repeating: 0, count: cells)
        used = 0
        for card in cards {
            let p0 = chroma ? alignedChromaPatch(card, template) : alignedPatch(card, template)
            guard let p = p0, let n = sparkleNormalise(p) else { continue }
            for i in 0..<cells { acc[i] += n[i] }
            used += 1
        }
        guard used > 0, let next = sparkleNormalise(acc.map { $0 / CGFloat(used) }) else {
            die("pass \(pass) produced no template")
        }
        FileHandle.standardError.write(Data(
            "pass \(pass): agreement with previous \(String(format: "%.4f", Double(sparkleCorrelate(next, template))))\n".utf8))
        template = next
    }

    var swift = """
    // The foil sparkle's \(chroma ? "warm-cool" : "luma") template. GENERATED — do not hand-edit.
    //
    //     make cardkit
    //     ./bin/cardkit-probe --sparkle-fit scan/foil-corpus \\
    //         scan/hoard-scan/Sources/BorderKit/Sparkle\(chroma ? "Chroma" : "")TemplateData.swift\(chroma ? " --chroma" : "")
    //
    // \(SparkleTemplate.cols)x\(SparkleTemplate.rows) cells spanning
    // SparkleTemplate.spanU x spanV of the card, zero-mean and unit-norm, so
    // correlating a normalised patch against it is a plain dot product. See
    // Sparkle.swift for what it is and why it is shaped this way.

    import CoreGraphics

    /// How many foil captures were averaged into this. Recorded because the
    /// threshold beside it is only as good as the corpus behind both.
    public let \(chroma ? "sparkleChromaTemplateFittedFrom" : "sparkleTemplateFittedFrom") = \(used)

    public let \(chroma ? "sparkleChromaTemplate" : "sparkleTemplate"): [CGFloat] = [\n
    """
    for j in 0..<SparkleTemplate.rows {
        let row = (0..<SparkleTemplate.cols)
            .map { String(format: "%.6f", Double(template[j * SparkleTemplate.cols + $0])) }
            .joined(separator: ", ")
        swift += "    \(row),\n"
    }
    swift += "]\n"

    do { try swift.write(toFile: out, atomically: true, encoding: .utf8) }
    catch { die("could not write \(out): \(error)") }
    print("fitted from \(used) foils\(onlySession.map { " (session \($0) only)" } ?? "") -> \(out)")
    exit(0)
}

func sparkleScoreCorpus(dir: String, verbose: Bool, onlySession: String?) -> Never {
    let labels = readLabels("\(dir)/labels.tsv")
        .filter { onlySession == nil || $0.session == onlySession! }
    var byClass: [String: [(CGFloat, CorpusRow)]] = [:]
    var maxSamples = 0

    var chromaByClass: [String: [(CGFloat, CorpusRow)]] = [:]
    for row in labels {
        guard let card = CorpusCard(URL(fileURLWithPath: "\(dir)/cards/\(row.id).png")),
              let r = sparkleScan(card.sampler) else {
            print("\(row.id)\tNO READING")
            continue
        }
        let c = sparkleScan(card.chromaSampler, template: sparkleChromaTemplate)
        maxSamples = max(maxSamples, r.samples)
        let key = row.frame == "modern" ? "modern" : "retro \(row.finish)"
        byClass[key, default: []].append((r.score, row))
        chromaByClass[key, default: []].append((c?.score ?? 0, row))
        if verbose {
            print(String(format: "%-7@ %-14@ %-8@ %7.3f  du %+.4f dv %+.4f  mad %.3f  chroma %7.3f  cmad %.3f",
                         row.id as NSString, row.name.prefix(14) as NSString,
                         row.finish as NSString, Double(r.score),
                         Double(r.offsetU), Double(r.offsetV), Double(r.contrast),
                         Double(c?.score ?? 0), Double(c?.contrast ?? 0)))
        }
    }

    print("\nthreshold \(SparkleGate.accept), template from \(sparkleTemplateFittedFrom) foils")
    print(String(format: "%-16@ %4@ %8@ %8@ %8@ %10@",
                 "class" as NSString, "n" as NSString, "min" as NSString,
                 "median" as NSString, "max" as NSString, "accepted" as NSString))
    for key in ["retro foil", "retro nonfoil", "modern"] {
        guard let rows = byClass[key], !rows.isEmpty else { continue }
        let v = rows.map(\.0).sorted()
        let accepted = v.filter { $0 >= SparkleGate.accept }.count
        print(String(format: "%-16@ %4d %8.3f %8.3f %8.3f %10d",
                     key as NSString, v.count, Double(v[0]),
                     Double(v[v.count / 2]), Double(v[v.count - 1]), accepted))
    }

    print("\nwarm-cool channel, threshold \(SparkleGate.acceptChroma)")
    for key in ["retro foil", "retro nonfoil", "modern"] {
        guard let rows = chromaByClass[key], !rows.isEmpty else { continue }
        let v = rows.map(\.0).sorted()
        let accepted = v.filter { $0 >= SparkleGate.acceptChroma }.count
        print(String(format: "%-16@ %4d %8.3f %8.3f %8.3f %10d",
                     key as NSString, v.count, Double(v[0]),
                     Double(v[v.count / 2]), Double(v[v.count - 1]), accepted))
    }

    print("\neither channel (measured only — chroma does not vote)")
    for key in ["retro foil", "retro nonfoil", "modern"] {
        guard let lum = byClass[key], let chr = chromaByClass[key], !lum.isEmpty else { continue }
        let accepted = zip(lum, chr).filter {
            $0.0 >= SparkleGate.accept || $1.0 >= SparkleGate.acceptChroma
        }.count
        print(String(format: "%-16@ %4d %30@ %10d",
                     key as NSString, lum.count, "" as NSString, accepted))
    }

    for (key, rows) in byClass.sorted(by: { $0.key < $1.key }) {
        guard let chr = chromaByClass[key] else { continue }
        let paired = Array(zip(rows, chr))
        let wrong = key == "retro foil"
            ? paired.filter { $0.0.0 < SparkleGate.accept && $0.1.0 < SparkleGate.acceptChroma }
            : paired.filter { $0.0.0 >= SparkleGate.accept || $0.1.0 >= SparkleGate.acceptChroma }
        for (l, c) in wrong.sorted(by: { max($0.0.0, $0.1.0) > max($1.0.0, $1.1.0) }) {
            let what = key == "retro foil" ? "MISSED foil" : "FALSE POSITIVE"
            print(String(format: "  %@: %@ %@ (luma %.3f, chroma %.3f)", what as NSString,
                         l.1.id as NSString, l.1.name as NSString,
                         Double(l.0), Double(c.0)))
        }
    }

    print("\npixel samples per card: \(maxSamples) (ceiling \(SparkleGate.maxSamples))")
    if maxSamples > SparkleGate.maxSamples {
        print("OVER BUDGET")
        exit(3)
    }
    exit(0)
}

@available(macOS 15, *)
func anchorFit(manifest: String) async -> Never {
    let dir = URL(fileURLWithPath: manifest).deletingLastPathComponent()
    guard let text = try? String(contentsOfFile: manifest, encoding: .utf8) else {
        die("no manifest at \(manifest)")
    }
    print("era\tframe\tkind\tprefix\tleftU\tsetCode\tnumberSource\tname")
    for row in text.split(whereSeparator: \.isNewline).dropFirst() {
        let f = row.split(separator: "\t", omittingEmptySubsequences: false).map(String.init)
        guard f.count >= 6 else { continue }
        let (sid, era, name) = (f[0], f[1], f[3])
        let img = dir.appendingPathComponent("images/\(sid).png")
        guard let src = CGImageSourceCreateWithURL(img as CFURL, nil),
              let cg = CGImageSourceCreateImageAtIndex(src, 0, nil) else { continue }

        guard let fit = await measureAnchorOnFlatCard(cg) else { continue }
        let m = fit.anchor
        print("\(era)\t\(fit.year)\t\(m.kind)\t\(m.prefix)"
            + "\t\(String(format: "%.4f", Double(m.leftU)))"
            + "\t\(fit.setCode.isEmpty ? "-" : fit.setCode)\t\(fit.numberSource)\t\(name)")
    }
    exit(0)
}

func sparkleWhere(path: String) async -> Never {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { die("could not read image: \(path)") }
    let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
        .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
        .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up
    let upright = uprighted(cg, orientation)
    guard let located = locateCard(upright) else {
        die("no card located in \(path)")
    }
    let flat = located.image

    let wide = SparkleWindow(
        u: SparkleGate.searchU * 4, v: SparkleGate.searchV * 4,
        cellsU: SparkleGate.searchCellsU * 4, cellsV: SparkleGate.searchCellsV * 4)

    let fitted = sparkleInCard(flat)
    let found = sparkleInCard(flat, window: wide)
    let fittedScore: Double = fitted?.luma.map { Double($0.score) } ?? -9
    let fittedU: Double = fitted?.luma.map { Double($0.offsetU) } ?? 0
    let fittedV: Double = fitted?.luma.map { Double($0.offsetV) } ?? 0
    let wideScore: Double = found?.luma.map { Double($0.score) } ?? -9
    let wideU: Double = found?.luma.map { Double($0.offsetU) } ?? 0
    let wideV: Double = found?.luma.map { Double($0.offsetV) } ?? 0
    let outside: Bool = abs(wideU) > Double(SparkleGate.searchU)
        || abs(wideV) > Double(SparkleGate.searchV)
    var out: [String: Any] = [:]
    out["file"] = (path as NSString).lastPathComponent
    out["fittedScore"] = fittedScore
    out["fittedU"] = fittedU
    out["fittedV"] = fittedV
    out["wideScore"] = wideScore
    out["wideU"] = wideU
    out["wideV"] = wideV
    out["outside"] = outside
    out["fittedContrast"] = fitted?.luma.map { Double($0.contrast) } ?? -9
    out["wideContrast"] = found?.luma.map { Double($0.contrast) } ?? -9
    out["fittedChroma"] = fitted?.chroma.map { Double($0.score) } ?? -9
    out["fittedChromaContrast"] = fitted?.chroma.map { Double($0.contrast) } ?? -9
    if let d = try? JSONSerialization.data(withJSONObject: out),
       let s = String(data: d, encoding: .utf8) {
        print(s)
    }
    exit(0)
}

func sparkleChromaTrial(dir: String) -> Never {
    let labels = readLabels("\(dir)/labels.tsv").filter { $0.frame == "retro" }
    func load(_ row: CorpusRow) -> CorpusCard? {
        CorpusCard(URL(fileURLWithPath: "\(dir)/cards/\(row.id).png"))
    }
    let cells = SparkleTemplate.cols * SparkleTemplate.rows

    for (fitOn, scoreOn) in [("s1", "s2"), ("s2", "s1")] {
        let fitRows = labels.filter { $0.session == fitOn && $0.finish == "foil" }
        let cards = fitRows.compactMap(load)
        guard !cards.isEmpty else { continue }

        var acc = [CGFloat](repeating: 0, count: cells)
        var used = 0
        for c in cards {
            guard let p = sparklePatch(du: 0, dv: 0, step: 1, c.chromaSampler),
                  let n = sparkleNormalise(p) else { continue }
            for i in 0..<cells { acc[i] += n[i] }
            used += 1
        }
        guard used > 0, var template = sparkleNormalise(acc.map { $0 / CGFloat(used) })
        else { continue }
        for _ in 1...3 {
            acc = [CGFloat](repeating: 0, count: cells)
            used = 0
            for c in cards {
                guard let p = alignedChromaPatch(c, template),
                      let n = sparkleNormalise(p) else { continue }
                for i in 0..<cells { acc[i] += n[i] }
                used += 1
            }
            guard used > 0, let next = sparkleNormalise(acc.map { $0 / CGFloat(used) })
            else { break }
            template = next
        }

        var byClass: [String: [CGFloat]] = [:]
        var perCard: [(CorpusRow, CGFloat, CGFloat)] = []
        for row in labels where row.session == scoreOn {
            guard let c = load(row),
                  let r = sparkleScanTemplate(c.chromaSampler, template: template) else { continue }
            byClass[row.finish, default: []].append(r.score)
            let l = sparkleScan(c.sampler)?.score ?? -9
            perCard.append((row, l, r.score))
        }
        for (row, l, ch) in perCard.sorted(by: { $0.0.finish + $0.0.name < $1.0.finish + $1.0.name }) {
            print(String(format: "    %-8@ %-22@ luma %6.3f  chroma %6.3f",
                         row.finish as NSString, row.name.prefix(22) as NSString,
                         Double(l), Double(ch)))
        }
        print("\nchroma template fitted on \(fitOn) (\(cards.count) foils), scored on \(scoreOn):")
        for k in ["foil", "nonfoil"] {
            guard let v = byClass[k]?.sorted(), !v.isEmpty else { continue }
            print(String(format: "  %-8@ n=%2d  min %6.3f  median %6.3f  max %6.3f",
                         k as NSString, v.count, Double(v[0]),
                         Double(v[v.count / 2]), Double(v[v.count - 1])))
        }
        if let f = byClass["foil"]?.sorted(), let n = byClass["nonfoil"]?.sorted(),
           !f.isEmpty, !n.isEmpty {
            let clean = f.filter { $0 > n[n.count - 1] }.count
            print("  foils above the highest nonfoil (\(String(format: "%.3f", Double(n[n.count - 1])))): \(clean) of \(f.count)")
        }
    }
    exit(0)
}

func alignedChromaPatch(_ card: CorpusCard, _ template: [CGFloat]) -> [CGFloat]? {
    let sample = card.chromaSampler
    let cellU = SparkleGate.searchU / CGFloat(SparkleGate.searchCellsU)
    let cellV = SparkleGate.searchV / CGFloat(SparkleGate.searchCellsV)
    var best = -CGFloat.infinity
    var bestI = 0, bestJ = 0
    for i in -SparkleGate.searchCellsU...SparkleGate.searchCellsU {
        for j in -SparkleGate.searchCellsV...SparkleGate.searchCellsV {
            guard let p = sparklePatch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV,
                                       step: 1, sample),
                  let n = sparkleNormalise(p) else { continue }
            let s = sparkleCorrelate(n, template)
            if s > best { best = s; bestI = i; bestJ = j }
        }
    }
    guard best > -CGFloat.infinity else { return nil }
    return sparklePatch(du: CGFloat(bestI) * cellU, dv: CGFloat(bestJ) * cellV, step: 1, sample)
}

func sparkleScanTemplate(_ sample: @escaping CardSampler,
                         template: [CGFloat]) -> (score: CGFloat, contrast: CGFloat)? {
    let cellU = SparkleGate.searchU / CGFloat(SparkleGate.searchCellsU)
    let cellV = SparkleGate.searchV / CGFloat(SparkleGate.searchCellsV)
    var best = -CGFloat.infinity
    var bestPatch: [CGFloat]? = nil
    for i in -SparkleGate.searchCellsU...SparkleGate.searchCellsU {
        for j in -SparkleGate.searchCellsV...SparkleGate.searchCellsV {
            guard let p = sparklePatch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV,
                                       step: 1, sample),
                  let n = sparkleNormalise(p) else { continue }
            let s = sparkleCorrelate(n, template)
            if s > best { best = s; bestPatch = p }
        }
    }
    guard best > -CGFloat.infinity, let p = bestPatch else { return nil }
    let mean = p.reduce(0, +) / CGFloat(p.count)
    let spread = p.reduce(0) { $0 + abs($1 - mean) } / CGFloat(p.count)
    return (best, spread)
}

@available(macOS 15, *)
func sparkleControl(path: String) async -> Never {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { die("could not read image: \(path)") }
    let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
        .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
        .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up
    let upright = uprighted(cg, orientation)
    guard let located = locateCard(upright) else {
        die("no card located in \(path)")
    }
    let flat = located.image

    func fields(_ v: SparkleVerdict?, into out: inout [String: Any], prefix: String) {
        out[prefix + "Score"] = v?.luma.map { Double($0.score) } ?? -9
        out[prefix + "Contrast"] = v?.luma.map { Double($0.contrast) } ?? -9
        out[prefix + "Chroma"] = v?.chroma.map { Double($0.score) } ?? -9
        out[prefix + "ChromaContrast"] = v?.chroma.map { Double($0.contrast) } ?? -9
    }

    var out: [String: Any] = ["file": (path as NSString).lastPathComponent]
    fields(sparkleInCard(flat), into: &out, prefix: "target")
    fields(sparkleInCard(flat, anchorShiftU: 0.10), into: &out, prefix: "controlR")
    fields(sparkleInCard(flat, anchorShiftU: -0.10), into: &out, prefix: "controlL")
    if let d = try? JSONSerialization.data(withJSONObject: out),
       let s = String(data: d, encoding: .utf8) {
        print(s)
    }
    exit(0)
}

@available(macOS 15, *)
func creditAnchor(path: String) async -> Never {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { die("could not read image: \(path)") }
    let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
        .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
        .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up
    let upright = uprighted(cg, orientation)
    guard let located = locateCard(upright) else { die("no card located in \(path)") }
    let flat = located.image

    var out: [String: Any] = ["file": (path as NSString).lastPathComponent,
                              "aspect": Double(flat.width) / Double(flat.height)]

    if let band = cropCard(flat, CardGeometry.band) {
        let lines = await recognizeLines(band, correctLanguage: false)
        func squash(_ s: String) -> String {
            String(s.lowercased().filter { $0.isLetter })
        }
        func cardBox(_ b: CGRect) -> [String: Double] {
            ["u0": Double(b.minX), "u1": Double(b.maxX),
             "vMid": Double(0.82 + (1 - b.midY) * 0.18),
             "vTop": Double(0.82 + (1 - b.maxY) * 0.18)]
        }
        if let credit = lines.first(where: { squash($0.text).contains("llu")
            || squash($0.text).hasPrefix("lus") || squash($0.text).hasPrefix("flu") }) {
            out["credit"] = cardBox(credit.box)
            out["creditText"] = credit.text
        }
        if let company = lines.first(where: {
            let s = squash($0.text)
            return s.contains("wizard") || s.contains("coast") || $0.text.contains("©")
        }) {
            out["company"] = cardBox(company.box)
            out["companyText"] = company.text
        }
    }

    let wide = SparkleWindow(
        u: SparkleGate.searchU * 4, v: SparkleGate.searchV * 4,
        cellsU: SparkleGate.searchCellsU * 4, cellsV: SparkleGate.searchCellsV * 4)
    if let fitted = sparkleInCard(flat)?.luma {
        out["fittedScore"] = Double(fitted.score)
        out["fittedU"] = Double(fitted.offsetU); out["fittedV"] = Double(fitted.offsetV)
        out["fittedContrast"] = Double(fitted.contrast)
    }
    if let wideR = sparkleInCard(flat, window: wide)?.luma {
        out["wideScore"] = Double(wideR.score)
        out["wideU"] = Double(wideR.offsetU); out["wideV"] = Double(wideR.offsetV)
    }
    if let company = out["company"] as? [String: Double], let vMid = company["vMid"] {
        let shiftV = CGFloat(vMid - 0.0671) - CardLayout.sparkleV
        if let anchored = sparkleInCard(flat, anchorShiftV: shiftV)?.luma {
            out["anchoredScore"] = Double(anchored.score)
            out["anchoredU"] = Double(anchored.offsetU)
            out["anchoredV"] = Double(anchored.offsetV)
            out["anchoredContrast"] = Double(anchored.contrast)
            out["anchorShiftV"] = Double(shiftV)
        }
    }
    if let d = try? JSONSerialization.data(withJSONObject: out),
       let s = String(data: d, encoding: .utf8) {
        print(s)
    }
    exit(0)
}

struct FlatCard {
    let width: Int, height: Int
    private let data: [UInt8]

    init?(_ cg: CGImage) {
        let w = cg.width, h = cg.height
        guard w > 0, h > 0 else { return nil }
        var buf = [UInt8](repeating: 0, count: w * h * 4)
        let ok = buf.withUnsafeMutableBytes { raw -> Bool in
            guard let ctx = CGContext(
                data: raw.baseAddress, width: w, height: h, bitsPerComponent: 8,
                bytesPerRow: w * 4, space: CGColorSpaceCreateDeviceRGB(),
                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
            return true
        }
        guard ok else { return nil }
        width = w; height = h; data = buf
    }

    func luma(_ u: CGFloat, _ v: CGFloat) -> CGFloat? {
        let x = Int((u * CGFloat(width)).rounded(.down))
        let y = Int((v * CGFloat(height)).rounded(.down))
        guard x >= 0, x < width, y >= 0, y < height else { return nil }
        let o = (y * width + x) * 4
        let r = CGFloat(data[o]) / 255, g = CGFloat(data[o + 1]) / 255,
            b = CGFloat(data[o + 2]) / 255
        return 0.2126 * r + 0.7152 * g + 0.0722 * b
    }
}

@available(macOS 15, *)
func sparkleShape(path: String) async -> Never {
    guard let src = CGImageSourceCreateWithURL(URL(fileURLWithPath: path) as CFURL, nil),
          let cg = CGImageSourceCreateImageAtIndex(src, 0, nil)
    else { die("could not read image: \(path)") }
    let orientation = CGImageSourceCopyPropertiesAtIndex(src, 0, nil)
        .flatMap { ($0 as NSDictionary)[kCGImagePropertyOrientation] as? UInt32 }
        .flatMap { CGImagePropertyOrientation(rawValue: $0) } ?? .up
    let upright = uprighted(cg, orientation)
    guard let located = locateCard(upright) else { die("no card located in \(path)") }
    guard let flat = FlatCard(located.image) else { die("no pixels in \(path)") }
    let sample: CardSampler = { u, v in flat.luma(u, v) }

    let cellU = SparkleGate.searchU / CGFloat(SparkleGate.searchCellsU)
    let cellV = SparkleGate.searchV / CGFloat(SparkleGate.searchCellsV)
    var best = -CGFloat.infinity
    var bestP: [CGFloat]? = nil
    for i in -SparkleGate.searchCellsU...SparkleGate.searchCellsU {
        for j in -SparkleGate.searchCellsV...SparkleGate.searchCellsV {
            guard let p = sparklePatch(du: CGFloat(i) * cellU, dv: CGFloat(j) * cellV,
                                       step: 1, sample),
                  let n = sparkleNormalise(p) else { continue }
            let s = sparkleCorrelate(n, sparkleTemplate)
            if s > best { best = s; bestP = p }
        }
    }
    var out: [String: Any] = ["file": (path as NSString).lastPathComponent]
    if let p = bestP {
        out["score"] = Double(best)
        let sorted = p.sorted()
        let median = sorted[p.count / 2]
        out["contrast"] = Double(medianAbsDev(p))
        let binP = p.map { $0 > median }
        let binT = sparkleTemplate.map { $0 > 0 }
        var agree = 0, interB = 0, unionB = 0
        for (a, b) in zip(binP, binT) {
            if a == b { agree += 1 }
            if a && b { interB += 1 }
            if a || b { unionB += 1 }
        }
        out["shapeAgree"] = Double(agree) / Double(p.count)
        out["shapeIoU"] = unionB > 0 ? Double(interB) / Double(unionB) : 0
        let cols = SparkleTemplate.cols, rowsN = SparkleTemplate.rows
        var rowMeans = [CGFloat](repeating: 0, count: rowsN)
        for j in 0..<rowsN {
            var sum: CGFloat = 0
            for i in 0..<cols { sum += p[j * cols + i] }
            rowMeans[j] = sum / CGFloat(cols)
        }
        let mean = rowMeans.reduce(0, +) / CGFloat(rowsN)
        let idxMean = CGFloat(rowsN - 1) / 2
        var num: CGFloat = 0, dR: CGFloat = 0, dI: CGFloat = 0
        for j in 0..<rowsN {
            let a = rowMeans[j] - mean, b = CGFloat(j) - idxMean
            num += a * b; dR += a * a; dI += b * b
        }
        let denom = (dR * dI).squareRoot()
        out["rowRamp"] = denom > 0 ? Double(num / denom) : 0
        let mid = rowsN / 3
        let midMean = rowMeans[mid..<(rowsN - mid)].reduce(0, +) / CGFloat(rowsN - 2 * mid)
        out["centerBump"] = Double(midMean - mean)
    }
    if let d = try? JSONSerialization.data(withJSONObject: out),
       let s = String(data: d, encoding: .utf8) {
        print(s)
    }
    exit(0)
}

func medianAbsDev(_ xs: [CGFloat]) -> CGFloat {
    func median(_ v: [CGFloat]) -> CGFloat {
        let s = v.sorted()
        return s.isEmpty ? 0 : s[s.count / 2]
    }
    let m = median(xs)
    return median(xs.map { abs($0 - m) })
}
